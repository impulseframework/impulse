# access

Go library for role-based access control (RBAC) with domain-partitioned permission management. The release's default roles travel with the release, in a role file embedded in the application binary; custom roles and role memberships live in typed tables owned by this library. Permission checks are answered by an immutable in-memory snapshot compiled from both — lock-free and allocation-free while the snapshot is fresh, and read again from the store, before the check answers, once it is older than its bound or a change was announced.

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

- **Scope**: Where a request is — the global partition (`accesstypes.GlobalScope()`) or one tenant domain (`accesstypes.DomainScope(domain)`). Permission checks, permission digests and the foothold question take a `Scope`, because a request is in exactly one partition. Global-ness is structural: no domain string means "global", so any tenant name is legal data (a tenant literally named "global" is an ordinary tenant). Scopes are opaque labels to access — the application owns its tenant list, and checks fail closed on unknown tenants. A scope that names nothing (the zero `accesstypes.Scope`, which `DomainScope` of an empty tenant also is) is refused with `accesstypes.ErrNoScope`: the code that asked made a mistake.
- **PolicyScope**: Where policy is held — a role membership, a custom role and its grants live in the global partition (`accesstypes.GlobalPolicyScope()`), in one tenant domain (`accesstypes.DomainPolicyScope(domain)`), or in every tenant domain (`accesstypes.EveryDomainPolicyScope()`). The policy store and the user manager take a `PolicyScope`. A membership held in every domain reaches every tenant, including a tenant the store holds no other row in, so a tenant created at run time needs nothing written for its every-domain members. Every domain is not a place a request can be in, so nothing converts a `PolicyScope` into a `Scope`, and passing a `PolicyScope` to a check fails to compile. `Scope.PolicyScope()` converts the other way, and `PolicyScope.Covers(scope)` answers whether policy held somewhere applies in a partition.
- **User**: Individual with assigned roles
- **Role**: Named collection of permissions, of one of two kinds. A default role is declared in the release's role file (see Default Roles): a global default is held in the global partition, a domain default in every tenant domain, and the store holds no row for either. A custom role is created at run time through the user manager and held where it was created; its identity is `(policy scope, role)`.
- **Permission**: Action that can be performed (List, Read, Create, Update, Delete, ...)
- **Resource**: What a permission applies to. A resource name has at most one dot: `employees` is a parent resource, `employees.name` is one field on it, and `employees.*` grants all fields by implication (covering fields that don't exist yet), except that `Update` on all fields is not `Update` on a field the release marks immutable. An endpoint grant (`employees`) gives no field visibility, and field grants don't grant the endpoint — that separation is the point of field-level control.

## Policy Stores

Each store owns six tables named `{Prefix}{Store}{Roles|UserRoles|RoleGrants|Groups|GroupMembers|GroupRoles}` — defaults yield `AccessRoles`, `AccessUserRoles`, `AccessRoleGrants`, `AccessGroups`, `AccessGroupMembers`, `AccessGroupRoles`. Applications with several independent permission stores in one database give each a store name (`WithStore("AdminPortal")` → `AccessAdminPortalRoles`, ...); separate tables make cross-store leakage structurally impossible.

The tables hold what is written at run time: custom roles, their grants, role memberships, and the groups of people with their members and the roles they hold. The release's default roles have no rows. Each row records where it is held in its own columns, never in a distinguished domain value: `Kind` is `global`, `domain` or `every` and is checked in the database, `DomainId` carries the tenant's id on a `domain` row and is empty otherwise, and `Axis` is the empty string on every row today. Any tenant name is therefore ordinary data.

The `access.Store` interface the two subpackages implement is sealed: only this module's stores satisfy it. The stores are thin; validation, change signaling and snapshot compilation live in the access package, one code path for every store. Both stores keep the same contract:

- `Roles` holds custom roles only, each with the description it was created with.
- `UserRoles` stands alone. A membership names its role by name alone, since a default role has no row to reference. `InsertUserRole` writes by role name and is how a default role is given. `InsertCustomUserRole` gives a custom role: it reads the role's row (the scope's own, or every domain's for one domain) in the transaction that writes the membership and writes nothing where the row is not there, so a role deleted at that moment is not given. An index led by the user (`{Prefix}{Store}UserRolesByUserID`) serves `ListUserMemberships`, which lists every membership a user holds wherever it is held.
- `RoleGrants` belongs to its custom role: interleaved in `Roles` on Spanner and a foreign key on PostgreSQL, both with cascading delete. A grant needs its role's row, and deleting a custom role deletes its grants.
- `DeleteRole` refuses while memberships held in that scope that have not ended still name the role, checked in the same transaction as the delete, and takes the ended ones with the role. A custom role given at the same moment takes its turn with the delete, by Spanner's serializable transactions and on PostgreSQL by the role's row, which the giver locks shared while it writes and the delete locks before it looks for who holds the role: either the delete sees the new row and refuses, or the giver finds the role gone and writes nothing. A role held in every domain is also refused while a membership or a group's role held in one domain names it and that domain has no custom role of the name itself, and the ended ones of those go with the role.
- A membership, a place in a group and a group's role each carry the period they count in, in two nullable columns, `StartsAt` and `EndsAt` (see Periods). An insert gives the row its period: it writes the row where the key is free, and where it is taken it replaces the period and leaves the rest of the row as it was. A membership carries the reason it was given for too, in a nullable column `Reason` (see What is held and why): its insert writes the reason with the period and replaces both where the key is taken, so the reason kept is the one the role was last given with. No row is written when a period begins or ends. The listings of what a person or a group holds return the rows that count at the database's present; `ReadPolicy` returns every row with its period.
- `Groups` holds the groups of people, each under an id the access package generates, with where it is kept (`Kind` is `global` or `domain`, checked in the database: no group is kept in every domain), its name and its description. A unique index (`{Prefix}{Store}GroupsByName`) holds a name to one group where the group is kept.
- `GroupMembers` holds the people in each group and `GroupRoles` the roles each group holds, a role named by name alone as a membership names it (`InsertCustomGroupRole` reads a custom role's row as `InsertCustomUserRole` does). Both reference their group (a foreign key in both stores), so `DeleteGroup` refuses a group that still has a member or holds a role that has not ended, checked in the same transaction as the delete, takes the ended ones with the group, and the database stands behind the check. A grant written to a role, or a person or a role given to a group, that was deleted at that moment is not written, and the caller is told the role or the group does not exist. An index led by the user (`{Prefix}{Store}GroupMembersByUserId`) serves the groups a person is in, and one led by where a role is held (`{Prefix}{Store}GroupRolesByScope`) serves the group roles held in one tenant.
- `ReadPolicy` reads the custom roles, their grants, the memberships, and the groups with their members and roles, with one consistent view of the store, for the snapshot to compile.
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

    "example.com/harbor/pkg/router" // the application's generated router
)

func main() {
    ctx := context.Background()

    pool, _ := pgxpool.New(ctx, "postgresql://user:pass@localhost/db")

    store, err := postgresstore.New(pool)
    if err != nil {
        log.Fatal(err)
    }

    // The collection is what the release declares, here Read on documents and
    // their fields: the application's generated router answers it. An
    // application that ships default roles adds
    // access.WithDefaultRoles(Roles(), Name); see Default Roles.
    client, err := access.New(store, router.Collection())
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

Permission checks are served from an immutable in-memory snapshot. A check that
finds the snapshot fresh is lock-free and allocation-free and does not touch the
database. Policy changes propagate between instances in near-realtime through a
change signal the application supplies (see Change signal below), and the
heartbeat interval (default 1m) bounds the staleness whatever happens to the
signal. Writes made through this client are visible to its own checks at once.

**Freshness is the check's own.** A check reads the policy store before it
answers in three cases: no snapshot is loaded yet; the snapshot's store read
began longer ago than the heartbeat interval; or a change was announced, by a
policy write on this instance or a hint from another, and no store read that
began after the announcement has landed. One read serves every check waiting at
that moment, so a burst of requests costs one read and not one each, and the
snapshot is swapped only when the content changed. A hint that arrives while a
read is already in flight is not cleared by that read: the next check reads
once more.

**The background loop is an accelerator.** It re-reads the store on every
heartbeat tick and on every hint, ahead of the checks, so on an instance that is
serving a check seldom finds the snapshot due and seldom reads. Nothing depends
on it. On a platform that gives a process CPU only while a request is in flight
(Cloud Run's default) the loop does not run between requests, and the first
check after an idle spell still answers from a store read no older than the
heartbeat interval, because the check makes that read itself. An instance can
record a hint only once it has CPU to receive it; one that sat idle is covered
by the interval until its listener has run.

**No read runs on the caller's context.** The engine reads the store under its
own context, with its own timeout (ten seconds), whoever asked. A check made
inside a database transaction is therefore safe: the Spanner client refuses a
second read under a transaction's context, and the engine's read is not under
it. A caller that goes away does not cancel a read other checks are waiting on.
A store that does not answer holds a check for less than twice the timeout: the
read in flight when the check came, and at most one after it.

**A read that fails** is reported to the reload-error hook and the last good
snapshot answers. The store is then left alone for one second: the checks that
come in that pause are answered from the last good snapshot without a read, so a
store that is down is read, and its failure reported, once a pause and not once
a check; a hint from another instance that arrives meanwhile stays recorded, and
the first check after the pause reads. A policy write on this instance ends the
pause, since a store that has just accepted a write is not down: the next check
reads it, and a read that fails after the write begins a pause of its own. The
pause counts from the moment the read's time ran out when the read is found
failed later than that, so on an instance that lost its CPU with a reload in
flight, and sat idle past that reload's timeout and the pause after it, the
first check reads the store itself. Only the first load has no snapshot to fall
back on: its failure is the check's error.

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
    // What the release declares: the generated router's collection. Every
    // grant's names are held to it, by one rule: a load skips and reports a
    // stored grant naming a permission, resource or field this release does
    // not have, instead of carrying it, and a write refuses one. A grant
    // that names a permission and no resource is held to the permission. A
    // client is not built without it.
    router.Collection(),
    // The release's default roles: the role file embedded in the binary,
    // validated against the collection, with the name of the auth whose
    // roles they are (see Default Roles).
    access.WithDefaultRoles(Roles(), Name),
    // Recommended: propagate changes between instances in near-realtime over
    // the application's signal channel (see Change signal).
    access.WithChangeSignal(policySignal),
    // Optional: tune the heartbeat interval, the bound on staleness
    // (default 1m).
    access.WithHeartbeatInterval(30*time.Second),
    // Optional: replace the hook that receives reload/signal failures and
    // the findings a load reports. The default writes each one to the
    // standard log; while reloads fail, checks keep serving the last good
    // snapshot.
    access.WithReloadErrorHandler(func(err error) { alert(err) }),
)

// Readiness: block until the first policy snapshot has loaded.
if err := client.WaitReady(ctx); err != nil { ... }

// Shutdown: stop reloading (checks keep serving the last snapshot, and read
// the store no more).
defer client.Close()
```

A grant the running release cannot use — a condition that does not parse, a
condition on a scope-wide grant that no scope-wide check could settle, a
permission, resource or field the release's collection does not declare, or, on a role's grant on a
resource, anything else a write of the grant is refused for (below) — does not
stop the load: it is left out of
the snapshot and reported to the reload-error hook as a `*access.SkippedGrant`.
The permission it would grant is denied, the rest of the policy loads, and the
instance becomes ready. A scope-wide grant names a permission and nothing else,
and is held to that: one whose permission the collection does not list is
skipped and reported like the rest, so it gives nobody the permission, is in no
listing of what the role holds, and is no foothold in its scope. That is what
makes a stored grant safe to outlive the release that wrote it: after a
rollback, or after a release removes a field or a permission, the grant is
denied and reported, and everything else keeps working. The row stays in the
store until it is removed (`DeleteRolePermission`, `DeleteRoleGrant`), so a
later release that declares the name again carries the grant again: the
report is the notice to remove a grant nobody means any more. A row no write
could have made (a dot in its resource or its field column, or a field and no
resource) is skipped and reported whatever the collection declares. No removal
by name reaches such a row: `DeleteAllRolePermissions` removes it with the
role's other grants, and `DeleteRole` with the role.

A load reports two more findings the same way. A custom role whose name a
default role of the running release has, in the same kind of scope, is a
`*access.ShadowedRole`: the default wins, the custom role's grants are skipped,
and its members hold the default role's grants; rename or delete the custom
role to clear it. A stored grant under a default role's name is skipped and
reported the same way where the store holds no row for the role. Memberships naming a role that is neither a default role nor
a custom role in their scope are an `*access.OrphanedMembership`: the members
hold nothing from it, as happens when a release retires a default role and its
memberships remain. Each finding reaches the hook once per compiled snapshot,
and `CheckPolicy` returns the same findings to the deploy (see Default Roles).

A custom role's grant set changes through `ChangeRoleGrants`, one store write
for the removals and the additions together, so a reader never sees a role
with neither its old grant nor its new one.

By the same collection, the writes that give a custom role a grant
(`AddRoleGrant`, `AddRoleGrants`, `AddRolePermissionResources`, the additions
of `ChangeRoleGrants`, and `AddRolePermission` for a grant that names no
resource) refuse a grant the release cannot use by its name: a
permission the release does not have, a permission it does not declare on the
resource or the field, and an action no role is given. The refusal is a bad
request that says which and why (`List on Ledgers cannot be given to a
role, because this release declares no List on Ledgers. ...`), and nothing
of the call is written, the rows beside the refused one included. The load
and the writes go by one rule for a grant's names, so the grants a load skips
by name are the ones another release wrote or that were written straight to
the store, never ones this release was asked for and took.

A write of a grant on a resource is held to the rest of the rule the role
file's grants are held to as well, so a role made at run time holds nothing a
default role could not. The resource is of the role's scope: a global resource
for a role held globally, a domain's for a role held in one domain or in every
domain, since a grant of another scope sits in a partition the role's holders
never look in. `Update` is not given on what the release marks immutable: a
grant that names such a field is refused, and `Update` on all of a resource's
fields (`Ledgers.*`) does not reach it. And
the condition parses and reads only the attributes the resource declares and
the subject vocabulary the application declares, a field's condition being
held to its resource's attributes. Each is refused as a bad request in the
role file's own sentence (`role Auditor is a domain role but grants Execute on
CloseLedger, a global-scoped resource — a role's grants live at its declared
scope; move the grant to a global role`, `role Auditor: Read grant on Ledgers:
condition references "owner", which is not an attribute of Ledgers`), and
nothing of the call is written. The load holds a role's stored grant on a
resource to the same rule: one that breaks it, such as a grant of another scope
or a condition that reads an attribute the release does not declare, is skipped
and reported as a `*access.SkippedGrant` with the write's reason. So such a
grant is no foothold where its role is held, no check meets a condition it
cannot resolve, and its holder's other grants decide as they did. The store
still holds the row, so `RoleGrants` lists it, marked with the reason
(`RoleGrant.Skipped`): a page of a role's grants shows it as one the role does
not hold, with why. A caller that reads the grants table itself, as the
generated list of the custom roles' grants does, asks the same of the rows it
read (`SkippedStoredGrants`): the answer for each is the reason, or nothing for
a grant its role holds, and a row stored under the name of a role the release
defines is answered as left out too.

A removal is held to nothing: a grant the release no longer declares can
always be taken away. No client is without
the collection: `New` takes it as an argument and refuses a nil one, a nil
pointer of a collection's type included, so there is no client that checks no
name. `access.IsNilCollection` is that check, exported for a package that
takes the collection as a setting and refuses a missing one in its own words,
naming the setting: the generated auth packages ask it before they open
anything.

### Change signal

The engine announces on the change signal once for every call that writes
policy, as the call's last step and before it returns, and watches the signal
for the hints other instances announce. A call of the `UserManager` announces
once however many rows it writes: giving a role to thirty people is one
announcement, and a call that fails part-way announces what it wrote. Each row
is visible to this instance's own checks the moment it is stored. The engine
leaves nothing to publish once the call has returned, so an instance that gets
no CPU once it has answered (Cloud Run's default) holds no announcement of the
engine's back, and a write that has returned before `Close` (a migrate
command's seed) has handed its announcement to the channel before the
application closes it. The channel is to do the same and return once the signal
is sent: the resource package's live service sends one write for the signals of
a kind that overlap, and each of them waits for the write that covers it. The
announcement has two seconds: one that fails or runs out is reported to the
reload-error hook, once for the call, and never fails the write, and the other
instances then learn of the change at their heartbeat interval. So a channel
that is down costs a call two seconds, whatever the call wrote.

A hint is recorded the moment it arrives, before any reload is tried, and wakes
the background loop: from then the next check reads the policy store before it
answers, whether or not the loop got there first, so a change that was
announced waits for no loop on an instance that heard the announcement; the old
snapshot answers only while the store cannot be read (see A read that fails,
above). The signal carries no policy, so a lost hint costs latency, bounded by
the heartbeat interval, and a duplicated one costs a read. Watch errors reach
the reload-error hook too.

The application supplies the signal as an adapter over its one signal channel,
through `access.ChangeSignalFunc(announce, watch)`. `announce` publishes the
policy kind on the channel and returns when its context ends, since the call
that wrote waits for it. `watch` subscribes to the policy kind, hands every
signal to the `onChange` the engine passes, and blocks until its context ends;
that is the `Watch` contract, and it keeps the subscription open for the life of
the client, its stop running when `watch` returns. Whenever `watch` returns
early (a subscription that ended), the engine re-invokes it after a delay, so a
`watch` that returns on connection loss is enough.

The heartbeat interval is the bound. With or without a signal, no check is
answered from a snapshot whose store read began longer ago than the interval
(default 1m, `WithHeartbeatInterval`): the loop re-reads on every tick of it,
and a check that finds it passed re-reads before it answers. So cross-instance
staleness is bounded by that interval while the signal is broken or absent, on
an instance that is serving and on one that sat idle alike. Correctness never
depends on the signal, and never on the loop.

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

client, err := access.New(store, router.Collection(), access.WithChangeSignal(policySignal))
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
deduplicate by sorted-Resources equality.

A scope-wide grant has no row for a condition to see, so the only condition
one can carry is about the time of the request (`now < '2027-03-01T00:00:00Z'`).
No write gives a scope-wide grant a condition: `AddRolePermission` takes none,
and a role file's grant always names a resource. One that has a condition is a
row put straight in the store. At snapshot load, a scope-wide grant whose
condition no scope-wide check could settle is left out and reported as a
`*access.SkippedGrant`, like any other grant the release cannot use: one that
reads a row, one that compares the time to a subject value, which only the
database can read, and one whose time the check cannot read (an instant that is
not RFC 3339, a zone the timezone database does not know, a time of day or a
day name that is none). The whole condition is held, as a role file's is: one
such part leaves the row out even where another part would settle the answer
first. Such a row gives nobody the permission, and its holder's other grants
decide as they did. One whose condition reads only the
time is kept and settled at check time against the `Environment`. So
`CheckUser` and `CheckRole` answer `Granted` or `Denied`, never `Conditional`.
A scope-wide check whose `Environment` lacks what a condition reads is a check
error, never a guess: the condition reads `now` and the `Environment` carries
no instant, or it reads the local zone and the `Environment` carries none.

A role file's grant on a resource carries a condition (`Condition`), so
`CheckUserResources` and `CheckRoleResources` do answer `Conditional`. A
condition that reads `now` is settled against the instant the `Environment`
carries (`accesstypes.EnvironmentAt`, or `WithNow`), sampled once per request;
without one the check is an error.

The role checks are the same seams evaluated against a role's effective
grants — its own and, transitively, every role it inherits — with no member
involved: `CheckRole` and `CheckRoleResources` answer exactly what
`CheckUser` and `CheckUserResources` answer a user holding only that role.
They serve sessions that operate *as a role* (an administrator working a
partner portal under a role chosen for the session — the session library's
impersonated sessions) as well as policy introspection. A role has no
identity of its own: a `subject` term in a row condition is bound by the
resource layer to the session's effective identity at render time, and a
scope-wide grant whose condition needs a subject is left out when the policy
loads, as it is for a user. `RoleHasGrants` and `RolePermissionDigest` are the role twins of the
user foothold and digest questions.

```go
env := accesstypes.NewEnvironment()

decisions, err := client.CheckUserResources(ctx, env, user, scope, "Read", "documents", "documents.title", "images")
decision, err := client.CheckUser(ctx, env, user, scope, "ExportReports") // scope-wide

decisions, err = client.CheckRoleResources(ctx, env, role, scope, "Update", "documents")
decision, err = client.CheckRole(ctx, env, role, scope, "ExportReports")
```

`ExportReports` here is a permission no resource registers: the application
checks it for the whole of a scope and for nothing else. A release has such a
permission because the application declares it, with `@manualAddPermission` on
a constant of type `accesstypes.Permission` in its resources package, and the
generated collection then lists it with no resource:

```go
// ExportReports is checked for the whole of a tenant.
//
// @manualAddPermission
const ExportReports accesstypes.Permission = "ExportReports"
```

A scope-wide grant of a permission the collection does not list is refused
when it is written and skipped when the policy is loaded, so a check of a
permission the application never declared answers `Denied` for everyone.

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
tenant in your own guard — your application owns the tenant table. The one
scope the check path refuses is the zero `accesstypes.Scope`, with
`accesstypes.ErrNoScope`.

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

`UserRoles` reports what the user holds where each role is held, so a membership
held in every domain is one entry under `EveryDomainPolicyScope()`, never one per
tenant. It is the user's own memberships and the roles of every group the user is
in (see Groups), each role once in each place: it is what a sign-in decides a
person by, so a person who holds a role through a group alone is somebody.
`UserPermissions` reports what the user holds in each scope from the roles held
there and in every domain, default and custom roles alike.

### Groups

A group of people holds roles the way a person does, and a person in a group holds
the group's roles wherever the group holds them, until they leave the group or the
group loses the role.

```go
mgr := client.UserManager()

// A group is kept for the whole application or in one tenant domain. The id
// is generated; the name is one group's where the group is kept.
watch, err := mgr.AddGroup(ctx, accesstypes.DomainPolicyScope("tenant1"), "Night watch", "The people on the night shift.")
auditors, err := mgr.AddGroup(ctx, accesstypes.GlobalPolicyScope(), "Auditors", "Reads every tenant's books.")

mgr.AddGroupRoles(ctx, watch, accesstypes.DomainPolicyScope("tenant1"), "Editor")
mgr.AddGroupRoles(ctx, auditors, accesstypes.EveryDomainPolicyScope(), "Viewer")
mgr.AddGroupUsers(ctx, watch, "john.doe", "jane.roe")

// Emptied, a group is deleted.
mgr.DeleteGroupUsers(ctx, watch, "john.doe", "jane.roe")
mgr.DeleteGroupRoles(ctx, watch, accesstypes.DomainPolicyScope("tenant1"), "Editor")
mgr.DeleteGroup(ctx, watch)
```

- A check costs what it did: the snapshot folds each group's roles into its members when
  it compiles, so a person in a group is checked exactly as a person who holds the role
  themselves. A role held both ways is held once, and losing one way leaves the other
- A group kept in one tenant domain is managed there and holds roles in that domain
  alone: `AddGroupRoles` and `GroupRole` refuse a role anywhere else. A group kept for the
  whole application holds roles in the global partition, in one domain or in every domain
- No group holds the Super Administrator role, so a group is no way around the rule that
  only a super administrator makes another: the writes refuse it
- `DeleteGroup` refuses a group that still has a member or holds a role. Nobody loses a
  role because a group was deleted: each person is removed and each role taken by an act
  of its own
- `DeleteRole` refuses a custom role a group holds, as it refuses one a person holds: a
  role deleted from under a group would leave the group's row behind, and a role of the
  name made again would at once be held by everyone in the group. Take the role away
  from the group first
- `DeleteRole` of a role held in every tenant refuses while a person or a group holds the
  role in one tenant. A tenant with a custom role of the same name of its own does not
  stand in the way: its people hold that one
- `RoleUsers` lists who holds a role in a scope: the people who hold it by a membership
  of their own and the people who hold it through a group, each once. `RoleHolders` says
  how each holds it, their own membership, the groups, or both. Removing a person's own
  membership (`DeleteUserRoles`, `DeleteRoleUsers`) leaves what they hold through a
  group: that goes when they leave the group or the group loses the role
- A row the rules refuse (written past this package) gives nobody a role: the snapshot
  leaves it out and `CheckPolicy` reports it (`SkippedGroupRole`)
- `Groups`, `UserGroups`, `GroupUsers` and `GroupRoles` read what the store holds;
  `GroupRole`, with `NewGroupRoleKey` and `GroupMemberKey`, is for a caller that writes
  the rows in a transaction of its own and then calls `Controller.PolicyChanged`, as
  `Membership` is for a person's own role

### Periods

A role assignment may carry a period, a start and an end, and counts only inside it: a
role taken for a task goes away by itself, and nobody has to remember to remove it.

```go
mgr := client.UserManager()
tenant1 := accesstypes.DomainPolicyScope("tenant1")

// Until Friday noon, from now: no start is from the moment the role is given.
friday := time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC)
period, err := accesstypes.NewPeriod(nil, &friday)
mgr.AssignUserRole(ctx, tenant1, "john.doe", "Editor", period, "Covering the release on Friday")

// Assigning again replaces the period and the reason; with none, the role stands until it is removed.
mgr.AddUserRoles(ctx, tenant1, "john.doe", "Editor")

// A person's place in a group and a group's role take a period the same way.
mgr.AssignGroupUser(ctx, watch, "jane.roe", period)
mgr.AssignGroupRole(ctx, watch, tenant1, "Editor", period)
```

- Each end of a period is optional: no start is from the moment the assignment is given,
  and no end is until it is removed. The start is the first instant the assignment counts
  and the end the first it does not. A period that ends at or before its start is refused
- It holds at the moment, and nothing is written when a period begins or ends. A check
  reads the assignments that count at its instant, the Environment's `now`, which is the
  one a grant's condition folds with, and the engine's own clock where the Environment
  carries none. `UserRoles`, which a sign-in decides a person by, `RoleUsers`, `GroupUsers`,
  `GroupRoles` and `UserGroups` leave out an assignment outside its period while its row
  still stands, read at the database's present
- A check still costs what it did. The snapshot holds the assignments that count in a
  window of time, from the latest start or end before its instant until the earliest after
  it, and the first check past the window compiles the next one from the records it
  already holds, with no store read. A store whose assignments carry no period compiles
  one snapshot for all time
- A person holds a group's role while their place in the group and the group's role both
  count
- Changing a period is assigning again: `AssignUserRole`, `AssignGroupUser` and
  `AssignGroupRole` give the period handed, and the `Add` methods give none. Ending an
  assignment now is removing it
- The Super Administrator role takes no end, and no start either (`ValidatePeriod`):
  there is always a super administrator, and a period could leave none
- An assignment that has ended stands in nobody's way: `DeleteRole` and `DeleteGroup`
  take the ended ones with what they delete, and refuse one whose period has not begun
  as they refuse one that counts
- Nothing here removes an ended row on a schedule. The identity library removes a
  person's ended assignments at their next sign-in; until then the row counts for nothing
- A caller that writes an assignment's row in a transaction of its own asks
  `ValidatePeriod` before it writes, as it asks `Membership` or `GroupRole` for the key

### What is held and why

A person's role may carry the reason it was given for, and the user manager answers
three questions a page asks of what is held: where each of a person's roles comes from,
what each group holds, and who loses a role a group stops holding.

```go
// The reason is the last argument of AssignUserRole: one line, empty for none.
mgr.AssignUserRole(ctx, tenant1, "john.doe", "Editor", period, "Covering the release on Friday")

held, err := mgr.UserRolesHeld(ctx, "john.doe")       // []access.HeldRole: their own with period and reason, and through each group
groups, err := mgr.GroupContents(ctx, tenant1)          // []access.GroupContent: each group with its people counted and its roles
losers, err := mgr.GroupRoleLosers(ctx, watch, tenant1, "Editor") // who loses Editor in tenant1 when the group stops holding it
```

- The reason is one line of plain text of at most `MaxReason` characters, kept less the
  space around it; a line break or a character that cannot be shown is refused
  (`ValidateReason`, which a caller that writes the row in a transaction of its own asks
  before it writes). It is not policy: no check reads it, and `ReadPolicy` does not carry
  it
- Nothing edits a reason by itself. Assigning again replaces the period and the reason
  together, and the `Add` methods give neither, so a role given again with no reason
  carries none
- `UserRolesHeld` lists a role once for each way the person holds it: a membership of
  their own (`Group` is nil), with its period and its reason, and each group they are in
  that holds the role (`Group` names it). The person's own are the memberships that have
  not ended, one whose period has not begun among them; the ones through a group are the
  ones `UserRoles` counts now
- `GroupContents` counts what has not ended, an assignment whose period has not begun
  among it, which is what `DeleteGroup` refuses a group for: a group listed with no
  people and no roles is one `DeleteGroup` removes
- `GroupRoleLosers` lists the people in the group now who hold the role there through
  this group and in no other way. A person who also holds it themselves, through another
  group, or in every tenant when the scope is one tenant, keeps it and is not listed.
  Nobody is listed while the group does not give the role: its period has not begun or
  has ended, or the role is one its members do not hold through it

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

users, err := mgr.RoleUsers(ctx, tenant1, "Administrator")     // who holds it, themselves or through a group
holders, err := mgr.RoleHolders(ctx, tenant1, "Administrator") // []access.RoleHolder: each with how they hold it
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
with no exact match is an error. Only the case of ASCII letters is set aside: a
letter from outside ASCII matches only itself, so a name written with a
look-alike letter (the Kelvin sign for `k`, the long s for `s`) finds no role.
It is how a name from outside the application,
a directory's group (a Google group is an email address, so its name is
lowercase), finds the role the role file spelled. A custom role held in one
domain alone cannot be assigned in every domain.

The library keeps two roles in a scope from differing only in case: the role
file is refused at `New` when it declares such a pair, and `AddRole` refuses a
custom role whose name matches an existing role of the scope without regard to
case, so a resolution
is ambiguous only where a pair came about another way: a release that brings a
default role whose name differs only in case from a custom role a tenant already
holds. `ResolveRole` then answers an error that names the roles, until one of them
is removed. Two such names added at the same moment do not both land: the store
refuses the second in the insert's own transaction. A role added in every
domain is held to the custom roles of each single domain as well, since a name
asked in one domain is read against both. `AddRole` also holds a custom role's
name to the one grammar
every role name follows — letters and digits starting with a capital letter, such
as `Administrator` or `TenantAdministrator` (see Default Roles) — and refuses a
name outside it with the same words the role file is refused with.

A default role's name is taken: `AddRole` refuses it, and `DeleteRole` refuses
to delete a default role. `DeleteRole` also refuses while users hold the role
in that scope, and, for a role held in every domain, while a person or a group
holds it in one domain. A custom role held in every domain is changed where it is held,
through `EveryDomainPolicyScope()`, not through one domain. A role deleted at the
moment it is given is not given: the call that gives it answers that the role does not
exist, and what the same call gave before it stays given.

### Permission Management

```go
// Resource- and field-specific permissions
mgr.AddRolePermissionResources(ctx, tenant1, "Editor", "Read", "documents", "documents.*")
mgr.DeleteRolePermissionResources(ctx, tenant1, "Editor", "Read", "documents.*")

// A scope-wide permission (not tied to any resource) is granted through the
// base-name method — there is no resource value that means it. The
// permission is one the release has: one its collection lists, because a
// resource registers it or because the application declares it with
// @manualAddPermission on a constant of type accesstypes.Permission.
mgr.AddRolePermission(ctx, tenant1, "Administrator", "CreateUsers")
mgr.DeleteRolePermission(ctx, tenant1, "Administrator", "CreateUsers")

// What the role holds where a request is, from the snapshot, inheritance
// folded: the file's grants for a default role, the store's for a custom one.
permissions, err := mgr.RolePermissions(ctx, accesstypes.DomainScope("tenant1"), "Administrator")

// The role's resource grants, each with the condition it is granted under
// (access.RoleGrant). A stored row this release cannot use is listed too, with
// why in Skipped: the load left it out, so the role does not hold it.
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
takes one role file: a second `WithDefaultRoles` among its options is refused,
since an engine holds the roles of one auth. The file says whose roles it holds,
in its `auth` key, and `New` takes it only under that name: a file handed over
under another auth's name, a file with no `auth` handed over under a name, and a
file with one handed over under none are refused, so one auth's roles never open
another auth's engine. `New` parses the file and
validates it against the collection the release declares: a file that does not
parse, names a role outside the grammar, leaves a role without a description,
declares a role twice or at both scopes, grants what the collection does not
declare, or carries a condition the vocabulary refuses fails `New`, so a
release whose default roles are wrong does not start.

A collection may mark an action as one no role is given, as the generator marks the
action of the create-your-tenant route (`NoRole: true` in the generated collection).
Nothing asks for a grant of such an action: it is registered only so that a permission
mask can refuse it to a session that acts as someone. A role file that grants one is
refused, in a sentence that names the role and the action and says to remove the grant,
and a grant of one that the store already holds for a custom role is skipped at the load
and reported by `CheckPolicy`, as a grant on a name this release does not declare. The
collection says which actions are marked through two optional methods,
`IsNoRoleAction(action)` and `NoRoleActions()`, which the generated collection and
`UnionCollection` carry; a collection without them marks none.

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

Embed the role file in the auth's package, which gives it beside its name:

```go
// Package staff is the auth of the application's staff.
package staff

import (
    _ "embed"

    "github.com/impulseframework/impulse/access"
)

// Name is the auth's name.
const Name = "staff"

//go:embed roles.json
var roleFile access.RoleFile

// Roles returns the auth's role file.
func Roles() access.RoleFile {
    return roleFile
}
```

Hand it to `New`, which validates it against the collection it is given:

```go
client, err := access.New(store, router.Collection(),
    access.WithDefaultRoles(Roles(), Name),
    access.WithChangeSignal(policySignal), // see Change signal
)
if err != nil {
    return err // includes a role file that does not parse or does not validate
}
```

The collection is given once, to `New`: the role file validates against it, and
the names of every grant the store holds or a write adds are held to it, a
grant on a resource or a field and a grant of a permission for the whole of a
scope alike.

The second argument of `WithDefaultRoles`, `auth`, is the name of the auth whose roles the
file holds, as the auth's package names it (`Name` in a package `impulse add auth`
renders). It says whose subject sets the file is held to: see `access.UnreadSetWarning`
below. `ValidateRoles` takes the same name. The file carries the same name in its `auth`
key, and `New` and `ValidateRoles` refuse the two when they differ.

An application hands a role file over in the auth's own package, among the options of the
`access.New` call that opens the auth's engine, where `Roles()` and `Name` are the package's own
(`access.WithDefaultRoles(Roles(), Name)`): `impulse check` takes a hand-over written anywhere
else for a finding, since nothing there ties the file to the engine it opens.

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
the kind: `access.GrantWarning`, `access.RequiredFieldWarning`,
`access.ConcealingKeyWarning` and `access.UnreadSetWarning` come from the role file, and `*access.SkippedGrant`, `*access.ShadowedRole` and
`*access.OrphanedMembership` from the store.

`access.UnreadSetWarning` names a subject set the application declares
(`@subjectSet`) that no role reads. A set is read when a grant's condition
names it (`hangarId IN subject.hangarsOpen`), or when a set that is read builds
on it in its `who:` part (`GroupID IN subject.crewGroups`). Nobody holds
anything through an unread set, so the line says to write the grant that reads
it. It is never a refusal: a set may be declared one change before its first
grant, and the role-file test accepts the warning until then. The set of a
person's groups that the generator declares for an auth is not flagged, since
nobody wrote it.

A set describes the people of one auth: its user column holds that auth's
people, and the set is looked up by their ids. An engine that is told its auth's
name takes no grant that reads another auth's set, whether the grant is in its
role file, written to a custom role, or found in the store. An application with
more than one auth says whose people each set holds (`auth: staff` in the
`@subjectSet`, the auth's name as its package names it; generation refuses a set
that says none). A run knows the auths its own outlets declare and the auth
packages in the tree of its module (a package directly under a directory named
`auth` that constructs a session Auth, the rule `impulse check` finds an auth
package by), so the generate program of one site knows another site's auth in
the same module with nothing declared for it. Each auth hands its own name with
its role file:

```go
access.WithDefaultRoles(Roles(), Name)
warnings, err := access.ValidateRoles(collection, membersRoles, members.Name)
```

The check of an auth looks at that auth's sets and at the sets that name no
auth, and never at another auth's, so a set is never flagged as unread in the
role file of an auth whose roles could not read it. The name is the one
generation knows the auth by, the name of its package, and, where the
collection's generation knew any auth, a file handed over under a name it did
not know is refused with the name given and the names known
(`PermissionCollection.Auths`; the generated collection lists the application's
auths as its run read them, and a union every site's). Two sites that say one
set name describes two different auths' people are refused where the union is
built (`UnionCollection`), naming the set and both auths: a role file reads a
set by its name alone, so one of the two sets takes another name, or both name
the same auth. Where the collections of a union together know several auths, a
set that does not say whose people it describes is refused there too, naming the
set: the union is where an application's sites meet, and it holds the rule for
the auths one site's generation could not see. A grant whose condition reads a
set that describes another auth's people is refused, naming the grant, the set
and both auths: in the role file when it is loaded (`New`, `ValidateRoles`), and
on a custom role when the grant is written. One found in the store is left out
when the policy is loaded, and reported. The set is looked up by the other
auth's ids, so it says nothing about the people who hold the role, whichever way
the condition tests it. A set follows a person through sets of its own auth
alone: generation refuses a `who:` that tests a set labeled for another auth,
the set of a person's groups that the generator declares for another auth
included. That set is named after its auth (`subject.staffGroups` for the auth
`staff`) and names its auth without being told. An application with one auth
need not write `auth:`: every set is that auth's. Where the collection knows
several auths, an engine is told whose roles it holds: `New` refuses an empty
name and refuses to open with no role file, and `ValidateRoles` refuses an empty
name. Where it knows one auth or none, an empty name holds no grant to an auth's
sets, and its check looks at every set.

### Behavior

- A global default role is held in the global partition; a domain default role is held in every tenant domain, including a tenant created after the release started, with nothing written for it
- A membership names a default role by name alone: a global default is assigned in the global partition, a domain default in one domain or in every domain
- `Roles` and `ResolveRole` include the default roles of the scope's kind, `Roles` with the file's description, and `RolePermissions` and `RoleGrants` answer a default role's grants from the file
- `AddRole` refuses a default role's name, `DeleteRole` refuses a default role, and the grant methods refuse to change one; copy its grants into a custom role under another name to change them
- A role whose file entry has no grants exists and holds nothing until a grant is authored
- Validates resources, permissions, and conditions against the collection before the client starts, and refuses a grant on a resource whose scope is not its role's
- Prevents update permissions on immutable resources
- Warns, without rejecting, when a role holds a conditional Delete, Update, or targeted Execute on a row it can neither Read nor List, a Create on a resource without a field every create must be given, or a conditional List on a concealing field the resource sorts or filters by (see Warnings)
- A release that introduces a default role under a custom role's name shadows the custom role, and a release that retires a default role leaves its memberships holding nothing; both are reported, and the store is left as it is

### The Super Administrator Role

Beside the role file's roles, every client defines one role of the framework's own:
`SuperAdministrator` (`accesstypes.SuperAdministrator`), the way back in when everything
else is broken. It is in no role file and needs none; a client built without a role file
defines it too.

- The name is reserved in any case: a role file that defines a role of that name, global
  or domain, does not validate (`RoleFile.Parse`, `ValidateRoles` and `New` refuse it, so
  `impulse check` and the release's start do), and `AddRole` refuses it as a custom role
  anywhere
- It is held in the global partition alone: `AddUserRoles`, `AddRoleUsers` and `Membership`
  refuse a membership of it in one tenant or in every tenant
- `Roles` lists it among the global roles with the framework's description, and it holds
  nothing of its own unless the client is built with
  `access.WithSuperAdministratorMethods(methods...)`: the methods that give and take the
  auth's roles, which the auth's generated management names
  (`<auth>identity.SuperAdministratorMethods()`). The role then holds Execute on each,
  under no condition, whatever the role file grants anybody, so a super administrator
  gives and takes any role of the auth for any person. `New` refuses a method the
  release's collection does not declare at the global scope with Execute.
  `Client.SuperAdministratorMethods` answers what the client was built with, and the
  identity library refuses an engine built without the methods its auth names
- `Client.UserIsSuperAdministrator` and `UserChecker.SuperAdministrator` answer whether a
  person holds it, from the snapshot, by the one test of who is a super administrator
  (`accesstypes.RoleCollection.HoldsSuperAdministrator`): the question a session's reach
  into a tenant asks of its actor, and the sign-in policy's first rule asks of the person
  signing in. A super administrator's session enters every tenant whatever the tenant's
  sign-in settings say. The one other person a tenant's sign-in settings do not keep out
  is that tenant's own administrator, in that tenant alone (see A Tenant's
  Administrators)
- Who may hold it is the identity library's rule, not this one's, where the application
  gives the roles: only a holder makes or removes another, a holder's account disabled
  or enabled included, or changes how another signs in, and the last holder who can
  sign in keeps the role and the account. Giving the role ends nothing the person
  holds: their sessions, tokens and remembered browsers stand
- In an auth whose roles come from its directory, the directory decides who is a super
  administrator: the session library's role synchronization gives the role to a person
  the directory names for it and takes it from one it no longer names, the last
  holder's included, as it does every role. `UserManager.ResolveRole` finds the role in
  the global partition alone, so the synchronization writes it nowhere the store would
  refuse it, and nobody is seeded as one
- No group holds it (see Groups): it is given to one person at a time, so the rule on who
  makes another super administrator has no way around it
- It takes no end and no start (see Periods): `AssignUserRole` and `ValidatePeriod`
  refuse any period on it, since there is always a super administrator and a period could
  leave none

### A Tenant's Administrators

An application names the roles whose holders administer a tenant with
`access.WithTenantAdministratorRoles(roles...)`, and the engine answers who administers
which tenant. It is the question a tenant's sign-in settings ask before they keep a
person out: a tenant that admits its people through its directory only still admits its
own administrators with a password and a second factor, so a tenant whose directory has a
problem can be mended by the people who run it (the session library's
`signin.TenantDeclaration.AdmitsAdministrator` is the decision, and its README says what
the person signs in with).

- A person administers a tenant when they hold one of the named roles in the tenant
  itself, inside its period (see Periods): by a membership of their own in the tenant's
  domain, or through a group kept in that tenant, which is written in the tenant, its
  people and its roles alike
- A role held in every tenant does not count, and neither does one a group kept for the
  whole application holds in the tenant: both are how the application's own people are
  seated everywhere, and a tenant's sign-in settings shut those people out with everyone
  else
- `Client.UserAdministersTenant` and `UserChecker.AdministersTenant` answer for one person
  and one tenant from the snapshot, on every request, so the answer follows a role taken
  away or a period that ended at the next request. `UserManager.TenantsAdministered`
  answers every tenant a person administers from the store, which is what the sign-in
  decision reads. Both come to one statement of where a role must be held to count
- `Client.TenantAdministratorRoles` answers what the client was built with. A client built
  without the option answers that nobody administers any tenant, and a generated auth
  package passes the role file's tenant administrator roles (`AdministratorRoles()`)

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

The second warning is about a create that can never be completed. A create writes the
fields the caller's Create grants name and no others, and the commit refuses a row whose
NOT NULL column nothing filled. The generated collection names, for each resource, the
fields a create must be given by the client: the column takes no NULL, no column default
and no `default_create_fn` fills it, it is no key and no field the server owns, and a
grant names it for Create. A role that holds Create on a resource, and none of whose
Create grants there covers one of those fields, creates no row by itself: the page draws
the create form from the grant, the person fills in everything they may, and the commit
answers 409 with nothing they can act on.

```
Warning: role FlightLead: Create on Sorties is granted without launchedAt, which every create of Sorties must be given (the column takes no NULL and nothing fills it for the client): a create under this role alone is refused at commit, whatever the person submits. Grant Create on the field, give the column a default, or accept this warning where the same people hold another role that grants it.
```

The check reads every Create grant the role holds on the resource, whatever condition each
is under, so it says that no create can succeed, never that some cannot. It is per role,
like the first: two roles the same person holds can cover the field between them, which a
role file cannot say, so the line is a warning and the release starts.

The third warning is about cost, not disclosure. A sort or filter on a conditionally
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
config, err := staff.Roles().Parse()
if err != nil {
    return err
}
warnings, err := access.ValidateRoles(router.Collection(), config, staff.Name)
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
`New`: the registries merge, the subject namespace is the union of the sites'
declarations, and every other question about a resource is answered by the first
collection registering it. A resource several sites serve must be declared identically
in each; `UnionCollection` refuses collections that disagree on a shared resource's
permissions (fields included), scope, immutability, computed marking, or method target.
An action one site marks as an action no role is given is no role's in the union, and a
site that registers the same name for roles is refused: a name is one or the other in
every site. Collections that declare one subject set in two different ways are refused,
with both collections and the first thing they differ in named: a role file reads a set
by its name alone, so the name holds the same people in every site. So are collections
that know two auths whose names are one name in PascalCase (`crew_ops` and `crewOps`):
generation names an auth's tables and its set of a person's groups by that form. A
collection passed as nil is refused with a sentence that says which argument it was.

```go
collection, err := access.UnionCollection(consolerouter.Collection(), portalrouter.Collection())
if err != nil {
    return err
}

client, err := access.New(store, collection, access.WithDefaultRoles(Roles(), Name))
```

### JSON Configuration

`auth` is the name of the auth whose roles the file holds, as the auth's package names it; a
file handed over under no name leaves it out.

```json
{
  "auth": "staff",
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
`"fields": ["*"]` is all of the resource's fields, the ones a later release adds among
them: the grant a write to a custom role spells `Documents.*`. `Update` on all the fields
is not `Update` on a field the release marks immutable.

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
